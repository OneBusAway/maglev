import java.io.Serializable;
import java.lang.reflect.Field;
import java.util.*;
import org.onebusaway.gtfs.model.AgencyAndId;
import org.onebusaway.gtfs.model.Stop;
import org.onebusaway.transit_data_federation.bundle.services.EntityReplacementStrategy;
import org.onebusaway.transit_data_federation.bundle.tasks.EntityReplacementStrategyFactory;
import org.onebusaway.transit_data_federation.bundle.tasks.EntityReplacementStrategyImpl;

// Prints the stop replacements Java's EntityReplacementStrategyFactory builds
// from a consolidation file, one "absorbed<TAB>canonical" line per entry,
// with agency and id separated by "|" so whitespace in ids stays visible.
public class JavaParity {
  public static void main(String[] args) throws Exception {
    EntityReplacementStrategyFactory factory = new EntityReplacementStrategyFactory();
    Map<Class<?>, String> mappings = new HashMap<>();
    mappings.put(Stop.class, args[0]);
    factory.setEntityMappings(mappings);
    EntityReplacementStrategy strategy;
    try {
      strategy = factory.create();
    } catch (Exception e) {
      System.out.println("ERROR\t" + e);
      return;
    }
    Field field = EntityReplacementStrategyImpl.class.getDeclaredField("_entityReplacement");
    field.setAccessible(true);
    @SuppressWarnings("unchecked")
    Map<Class<?>, Map<Serializable, Serializable>> all =
        (Map<Class<?>, Map<Serializable, Serializable>>) field.get(strategy);
    Map<Serializable, Serializable> stops = all.getOrDefault(Stop.class, Map.of());
    List<String> lines = new ArrayList<>();
    for (Map.Entry<Serializable, Serializable> e : stops.entrySet()) {
      lines.add(format((AgencyAndId) e.getKey()) + "\t" + format((AgencyAndId) e.getValue()));
    }
    Collections.sort(lines);
    lines.forEach(System.out::println);
  }

  private static String format(AgencyAndId id) {
    return "[" + id.getAgencyId() + "|" + id.getId() + "]";
  }
}

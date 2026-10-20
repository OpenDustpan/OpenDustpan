

# UnitDetails

## oneOf schemas
* [OpenTofuDetails](OpenTofuDetails.md)
* [PulumiDetails](PulumiDetails.md)

## Example
```java
// Import classes:
import org.opendustpan.client.model.UnitDetails;
import org.opendustpan.client.model.OpenTofuDetails;
import org.opendustpan.client.model.PulumiDetails;

public class Example {
    public static void main(String[] args) {
        UnitDetails exampleUnitDetails = new UnitDetails();

        // create a new OpenTofuDetails
        OpenTofuDetails exampleOpenTofuDetails = new OpenTofuDetails();
        // set UnitDetails to OpenTofuDetails
        exampleUnitDetails.setActualInstance(exampleOpenTofuDetails);
        // to get back the OpenTofuDetails set earlier
        OpenTofuDetails testOpenTofuDetails = (OpenTofuDetails) exampleUnitDetails.getActualInstance();

        // create a new PulumiDetails
        PulumiDetails examplePulumiDetails = new PulumiDetails();
        // set UnitDetails to PulumiDetails
        exampleUnitDetails.setActualInstance(examplePulumiDetails);
        // to get back the PulumiDetails set earlier
        PulumiDetails testPulumiDetails = (PulumiDetails) exampleUnitDetails.getActualInstance();
    }
}
```


